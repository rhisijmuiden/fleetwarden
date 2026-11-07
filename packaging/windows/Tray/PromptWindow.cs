using System;
using System.ComponentModel;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Markup;
using System.Windows.Media;
using System.Windows.Media.Animation;
using System.Windows.Threading;

namespace FleetWarden.Tray
{
    // The pop-up at the bottom right of the screen. It cannot be closed
    // without a choice; when the countdown ends the agent goes ahead.
    public sealed class PromptWindow : Window
    {
        const string Layout = @"
<Border xmlns='http://schemas.microsoft.com/winfx/2006/xaml/presentation'
        xmlns:x='http://schemas.microsoft.com/winfx/2006/xaml'
        Margin='16' CornerRadius='12' Background='{DynamicResource Surface}' BorderBrush='{DynamicResource Line}' BorderThickness='1'
        TextElement.FontFamily='Segoe UI Variable Text, Segoe UI' TextElement.Foreground='{DynamicResource Fg}'>
  <Border.Effect>
    <DropShadowEffect BlurRadius='28' ShadowDepth='6' Direction='270' Opacity='0.28' Color='#000000'/>
  </Border.Effect>
  <Border.Resources>
    <Style x:Key='Btn' TargetType='Button'>
      <Setter Property='Cursor' Value='Hand'/>
      <Setter Property='Height' Value='34'/>
      <Setter Property='Padding' Value='14,0'/>
      <Setter Property='FontSize' Value='13'/>
      <Setter Property='FontWeight' Value='SemiBold'/>
      <Setter Property='BorderThickness' Value='1'/>
      <Setter Property='FocusVisualStyle' Value='{x:Null}'/>
      <Setter Property='Template'>
        <Setter.Value>
          <ControlTemplate TargetType='Button'>
            <Border x:Name='Bd' CornerRadius='8' Background='{TemplateBinding Background}' BorderBrush='{TemplateBinding BorderBrush}'
                    BorderThickness='{TemplateBinding BorderThickness}' Padding='{TemplateBinding Padding}'>
              <ContentPresenter HorizontalAlignment='Center' VerticalAlignment='Center'/>
            </Border>
            <ControlTemplate.Triggers>
              <Trigger Property='IsMouseOver' Value='True'><Setter TargetName='Bd' Property='Opacity' Value='0.88'/></Trigger>
              <Trigger Property='IsPressed' Value='True'><Setter TargetName='Bd' Property='Opacity' Value='0.75'/></Trigger>
              <Trigger Property='IsKeyboardFocused' Value='True'><Setter TargetName='Bd' Property='BorderBrush' Value='{DynamicResource Fg}'/></Trigger>
            </ControlTemplate.Triggers>
          </ControlTemplate>
        </Setter.Value>
      </Setter>
    </Style>
  </Border.Resources>
  <Grid>
    <Grid.RowDefinitions>
      <RowDefinition Height='Auto'/>
      <RowDefinition Height='Auto'/>
    </Grid.RowDefinitions>
    <Border Height='4' CornerRadius='11,11,0,0' Background='{DynamicResource Accent}'/>
    <StackPanel Grid.Row='1' Margin='20,14,20,18'>
      <StackPanel Orientation='Horizontal'>
        <Viewbox Width='22' Height='22'>
          <Canvas Width='64' Height='64'>
            <Path Fill='{DynamicResource Accent}' Data='M32,4 L8,13 L8,30 C8,45.5 18.2,56.6 32,60 C45.8,56.6 56,45.5 56,30 L56,13 Z'/>
            <Path Fill='{DynamicResource Inner}' Data='M32,10.5 L14,17.3 L14,30 C14,42 21.6,50.9 32,53.9 C42.4,50.9 50,42 50,30 L50,17.3 Z'/>
            <Path Stroke='{DynamicResource Dots}' StrokeThickness='2.2' StrokeStartLineCap='Round' StrokeEndLineCap='Round'
                  Data='M32,28.5 L32,33.5 M29,27 L24,34.5 M35,27 L40,34.5'/>
            <Ellipse Canvas.Left='27.5' Canvas.Top='19.5' Width='9' Height='9' Fill='#FFFFFF'/>
            <Ellipse Canvas.Left='18' Canvas.Top='34' Width='8' Height='8' Fill='{DynamicResource Dots}'/>
            <Ellipse Canvas.Left='38' Canvas.Top='34' Width='8' Height='8' Fill='{DynamicResource Dots}'/>
          </Canvas>
        </Viewbox>
        <TextBlock x:Name='Product' Margin='8,0,0,0' VerticalAlignment='Center' FontSize='12' Foreground='{DynamicResource Muted}'/>
      </StackPanel>
      <TextBlock x:Name='Heading' Margin='0,12,0,0' FontSize='16' FontWeight='SemiBold' TextWrapping='Wrap'/>
      <TextBlock x:Name='Body' Margin='0,6,0,0' FontSize='13' LineHeight='19' TextWrapping='Wrap' Foreground='{DynamicResource Muted}'/>
      <TextBlock x:Name='Countdown' Margin='0,14,0,0' FontSize='13' FontWeight='SemiBold'/>
      <Grid Margin='0,6,0,0' Height='4'>
        <Border CornerRadius='2' Background='{DynamicResource Track}'/>
        <Border x:Name='Bar' CornerRadius='2' Background='{DynamicResource Accent}' RenderTransformOrigin='0,0.5'>
          <Border.RenderTransform><ScaleTransform ScaleX='1'/></Border.RenderTransform>
        </Border>
      </Grid>
      <TextBlock x:Name='Remaining' Margin='0,10,0,0' FontSize='12' TextWrapping='Wrap' Foreground='{DynamicResource Muted}'/>
      <StackPanel Margin='0,16,0,0' Orientation='Horizontal' HorizontalAlignment='Right'>
        <Button x:Name='PostponeButton' Style='{StaticResource Btn}' Margin='0,0,8,0'
                Background='{DynamicResource Surface2}' BorderBrush='{DynamicResource Line}' Foreground='{DynamicResource Fg}'/>
        <Button x:Name='NowButton' Style='{StaticResource Btn}'
                Background='{DynamicResource Accent}' BorderBrush='{DynamicResource Accent}' Foreground='{DynamicResource AccentFg}'/>
      </StackPanel>
    </StackPanel>
  </Grid>
</Border>";

        public Prompt Prompt { get; }
        readonly Action<bool> answer;
        readonly DateTime deadline;
        readonly DispatcherTimer timer;
        readonly TextBlock countdown;
        readonly ScaleTransform barScale;
        bool answered;

        public PromptWindow(Prompt prompt, Action<bool> onAnswer, bool animate = true)
        {
            Prompt = prompt;
            answer = onAnswer;
            Title = Strings.Product(prompt);
            WindowStyle = WindowStyle.None;
            AllowsTransparency = true;
            Background = Brushes.Transparent;
            ResizeMode = ResizeMode.NoResize;
            Topmost = true;
            ShowInTaskbar = false;
            ShowActivated = false;
            SizeToContent = SizeToContent.Height;
            Width = 432; // 400 plus the shadow margin
            Theme.Apply(Resources, prompt.AccentColor);

            var root = (FrameworkElement)XamlReader.Parse(Layout);
            Content = root;
            T(root, "Product").Text = Strings.ManagedBy(prompt);
            T(root, "Heading").Text = Strings.Title(prompt);
            T(root, "Body").Text = Strings.Body(prompt);
            T(root, "Remaining").Text = Strings.PostponesLeft(prompt);
            countdown = T(root, "Countdown");
            barScale = (ScaleTransform)((Border)root.FindName("Bar")).RenderTransform;

            var now = (Button)root.FindName("NowButton");
            now.Content = Strings.Now(prompt);
            now.Click += (s, e) => Answer(false);
            var later = (Button)root.FindName("PostponeButton");
            later.Content = Strings.Postpone(prompt);
            later.Visibility = prompt.CanPostpone ? Visibility.Visible : Visibility.Collapsed;
            later.Click += (s, e) => Answer(true);

            deadline = DateTime.UtcNow.AddSeconds(Math.Max(prompt.CountdownSeconds, 1));
            timer = new DispatcherTimer { Interval = TimeSpan.FromMilliseconds(250) };
            timer.Tick += (s, e) => Tick();
            Tick();
            timer.Start();

            Loaded += (s, e) => Place(animate);
            SizeChanged += (s, e) => { if (IsLoaded) Place(false); };
        }

        static TextBlock T(FrameworkElement root, string name) => (TextBlock)root.FindName(name);

        void Tick()
        {
            var left = deadline - DateTime.UtcNow;
            if (left <= TimeSpan.Zero)
            {
                Answer(false); // nobody chose: go ahead
                return;
            }
            countdown.Text = Strings.Countdown(Prompt, TimeSpan.FromSeconds(Math.Ceiling(left.TotalSeconds)));
            barScale.ScaleX = Math.Max(0, Math.Min(1, left.TotalSeconds / Math.Max(Prompt.CountdownSeconds, 1)));
        }

        void Place(bool animate)
        {
            var area = SystemParameters.WorkArea;
            Left = area.Right - ActualWidth;
            var top = area.Bottom - ActualHeight;
            if (!animate)
            {
                Top = top;
                return;
            }
            Top = top + 24;
            Opacity = 0;
            var ease = new CubicEase { EasingMode = EasingMode.EaseOut };
            BeginAnimation(TopProperty, new DoubleAnimation(top, TimeSpan.FromMilliseconds(260)) { EasingFunction = ease, FillBehavior = FillBehavior.Stop });
            BeginAnimation(OpacityProperty, new DoubleAnimation(1, TimeSpan.FromMilliseconds(200)));
            Top = top;
        }

        void Answer(bool postpone)
        {
            if (answered) return;
            answered = true;
            timer.Stop();
            answer?.Invoke(postpone && Prompt.CanPostpone);
            Close();
        }

        // CloseQuietly closes the pop-up without an answer (the agent closed
        // the prompt, or the connection to the agent is gone).
        public void CloseQuietly()
        {
            answered = true;
            timer.Stop();
            Close();
        }

        protected override void OnClosing(CancelEventArgs e)
        {
            if (!answered) e.Cancel = true; // Alt+F4 is no choice
            base.OnClosing(e);
        }

        public void Flash() => Activate();
    }
}
