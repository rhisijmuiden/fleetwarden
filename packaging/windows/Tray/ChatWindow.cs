using System;
using System.ComponentModel;
using System.Globalization;
using System.Threading.Tasks;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Markup;
using System.Windows.Media;

namespace FleetWarden.Tray
{
    // ChatWindow is the chat with the IT department, at the bottom right of
    // the screen. The technician starts it; the user answers. Closing only
    // hides it: the next message of the technician shows it again.
    public sealed class ChatWindow : Window
    {
        const string Layout = @"
<Border xmlns='http://schemas.microsoft.com/winfx/2006/xaml/presentation'
        xmlns:x='http://schemas.microsoft.com/winfx/2006/xaml'
        Margin='16' CornerRadius='14' Background='{DynamicResource Surface}' BorderBrush='{DynamicResource Line}' BorderThickness='1'
        TextElement.FontFamily='Segoe UI Variable Text, Segoe UI' TextElement.Foreground='{DynamicResource Fg}' TextElement.FontSize='13'>
  <Border.Effect>
    <DropShadowEffect BlurRadius='24' ShadowDepth='4' Direction='270' Opacity='0.28' Color='#000000'/>
  </Border.Effect>
  <Border.Resources>
    <Style x:Key='Btn' TargetType='Button'>
      <Setter Property='Cursor' Value='Hand'/>
      <Setter Property='Height' Value='36'/>
      <Setter Property='Padding' Value='14,0'/>
      <Setter Property='FontWeight' Value='SemiBold'/>
      <Setter Property='Background' Value='{DynamicResource Accent}'/>
      <Setter Property='Foreground' Value='{DynamicResource AccentFg}'/>
      <Setter Property='BorderThickness' Value='0'/>
      <Setter Property='FocusVisualStyle' Value='{x:Null}'/>
      <Setter Property='Template'>
        <Setter.Value>
          <ControlTemplate TargetType='Button'>
            <Border x:Name='Bd' CornerRadius='8' Background='{TemplateBinding Background}' Padding='{TemplateBinding Padding}'>
              <ContentPresenter HorizontalAlignment='Center' VerticalAlignment='Center'/>
            </Border>
            <ControlTemplate.Triggers>
              <Trigger Property='IsMouseOver' Value='True'><Setter TargetName='Bd' Property='Opacity' Value='0.88'/></Trigger>
              <Trigger Property='IsPressed' Value='True'><Setter TargetName='Bd' Property='Opacity' Value='0.75'/></Trigger>
              <Trigger Property='IsEnabled' Value='False'><Setter TargetName='Bd' Property='Opacity' Value='0.45'/></Trigger>
            </ControlTemplate.Triggers>
          </ControlTemplate>
        </Setter.Value>
      </Setter>
    </Style>
    <Style x:Key='Close' TargetType='Button'>
      <Setter Property='Cursor' Value='Hand'/>
      <Setter Property='Width' Value='30'/>
      <Setter Property='Height' Value='30'/>
      <Setter Property='Foreground' Value='{DynamicResource AccentFg}'/>
      <Setter Property='FocusVisualStyle' Value='{x:Null}'/>
      <Setter Property='Template'>
        <Setter.Value>
          <ControlTemplate TargetType='Button'>
            <Border x:Name='Bd' CornerRadius='8' Background='Transparent'>
              <Path Data='M0,0 L10,10 M10,0 L0,10' Stroke='{TemplateBinding Foreground}' StrokeThickness='1.6' HorizontalAlignment='Center' VerticalAlignment='Center'/>
            </Border>
            <ControlTemplate.Triggers>
              <Trigger Property='IsMouseOver' Value='True'><Setter TargetName='Bd' Property='Background' Value='#22FFFFFF'/></Trigger>
            </ControlTemplate.Triggers>
          </ControlTemplate>
        </Setter.Value>
      </Setter>
    </Style>
    <Style x:Key='Box' TargetType='TextBox'>
      <Setter Property='Foreground' Value='{DynamicResource Fg}'/>
      <Setter Property='CaretBrush' Value='{DynamicResource Fg}'/>
      <Setter Property='Background' Value='{DynamicResource Surface}'/>
      <Setter Property='BorderBrush' Value='{DynamicResource Line}'/>
      <Setter Property='Padding' Value='8,8'/>
      <Setter Property='Template'>
        <Setter.Value>
          <ControlTemplate TargetType='TextBox'>
            <Border x:Name='Bd' CornerRadius='8' Background='{TemplateBinding Background}' BorderBrush='{TemplateBinding BorderBrush}' BorderThickness='1'>
              <ScrollViewer x:Name='PART_ContentHost' Margin='{TemplateBinding Padding}' VerticalScrollBarVisibility='Auto'/>
            </Border>
            <ControlTemplate.Triggers>
              <Trigger Property='IsKeyboardFocused' Value='True'><Setter TargetName='Bd' Property='BorderBrush' Value='{DynamicResource Accent}'/></Trigger>
            </ControlTemplate.Triggers>
          </ControlTemplate>
        </Setter.Value>
      </Setter>
    </Style>
  </Border.Resources>
  <Grid>
    <Grid.RowDefinitions>
      <RowDefinition Height='Auto'/>
      <RowDefinition Height='*'/>
      <RowDefinition Height='Auto'/>
    </Grid.RowDefinitions>
    <Border x:Name='Header' Grid.Row='0' CornerRadius='14,14,0,0' Background='{DynamicResource Accent}' Padding='16,12,8,12'>
      <DockPanel>
        <Button x:Name='CloseBtn' DockPanel.Dock='Right' Style='{StaticResource Close}' VerticalAlignment='Top'/>
        <StackPanel>
          <TextBlock x:Name='Title' FontSize='15' FontWeight='SemiBold' Foreground='{DynamicResource AccentFg}'/>
          <TextBlock x:Name='Sub' FontSize='12' Margin='0,2,0,0' Foreground='{DynamicResource AccentFg}' Opacity='0.85'/>
        </StackPanel>
      </DockPanel>
    </Border>
    <ScrollViewer x:Name='Scroll' Grid.Row='1' VerticalScrollBarVisibility='Auto' Padding='14,12' Background='{DynamicResource Surface2}'>
      <StackPanel x:Name='Lines'/>
    </ScrollViewer>
    <Border Grid.Row='2' BorderBrush='{DynamicResource Line}' BorderThickness='0,1,0,0' Padding='12'>
      <StackPanel>
        <TextBlock x:Name='Status' FontSize='12' Margin='2,0,0,8' Foreground='{DynamicResource Muted}' TextWrapping='Wrap' Visibility='Collapsed'/>
        <DockPanel x:Name='Compose'>
          <Button x:Name='Send' DockPanel.Dock='Right' Style='{StaticResource Btn}' Margin='8,0,0,0' VerticalAlignment='Bottom'/>
          <TextBox x:Name='Input' Style='{StaticResource Box}' AcceptsReturn='False' TextWrapping='Wrap' MaxLength='2000' MaxHeight='96' MinHeight='36'/>
        </DockPanel>
      </StackPanel>
    </Border>
  </Grid>
</Border>";

        readonly Func<string, Task<SelfServiceReply>> send;
        readonly FrameworkElement root;
        readonly StackPanel lines;
        readonly ScrollViewer scroll;
        readonly TextBox input;
        readonly Button sendBtn;
        readonly TextBlock title, sub, status;
        readonly FrameworkElement compose;
        long lastLine;
        bool quitting, sending;

        public ChatState State { get; private set; }

        public ChatWindow(Func<string, Task<SelfServiceReply>> onSend)
        {
            send = onSend;
            WindowStyle = WindowStyle.None;
            AllowsTransparency = true;
            Background = Brushes.Transparent;
            ResizeMode = ResizeMode.NoResize;
            ShowInTaskbar = true;
            Topmost = true;
            Width = 400;
            Height = 520;
            Theme.Apply(Resources, null);
            root = (FrameworkElement)XamlReader.Parse(Layout);
            Content = root;
            lines = (StackPanel)root.FindName("Lines");
            scroll = (ScrollViewer)root.FindName("Scroll");
            input = (TextBox)root.FindName("Input");
            sendBtn = (Button)root.FindName("Send");
            title = (TextBlock)root.FindName("Title");
            sub = (TextBlock)root.FindName("Sub");
            status = (TextBlock)root.FindName("Status");
            compose = (FrameworkElement)root.FindName("Compose");
            title.Text = SsText.ChatTitle;
            Title = SsText.ChatTitle;
            sendBtn.Content = SsText.ChatSend;
            System.Windows.Automation.AutomationProperties.SetName(input, SsText.ChatPlaceholder);
            ((Button)root.FindName("CloseBtn")).Click += (s, e) => Hide();
            ((FrameworkElement)root.FindName("Header")).MouseLeftButtonDown += (s, e) => { if (e.ButtonState == MouseButtonState.Pressed) DragMove(); };
            sendBtn.Click += async (s, e) => await SendInput();
            input.KeyDown += async (s, e) =>
            {
                if (e.Key == Key.Enter && (Keyboard.Modifiers & ModifierKeys.Shift) == 0)
                {
                    e.Handled = true;
                    await SendInput();
                }
            };
            input.TextChanged += (s, e) => sendBtn.IsEnabled = !sending && !string.IsNullOrWhiteSpace(input.Text) && State != null && State.Open;
            Loaded += (s, e) => Place();
        }

        void Place()
        {
            var area = SystemParameters.WorkArea;
            Left = area.Right - Width;
            Top = area.Bottom - Height;
        }

        // Apply shows a new state. It reports whether there is a new message
        // of the technician, so the caller can bring the window forward.
        public bool Apply(ChatState c)
        {
            var isNew = State == null || State.Id != c.Id;
            State = c;
            Theme.Apply(Resources, c.AccentColor);
            sub.Text = SsText.ChatWith(c.Operator, string.IsNullOrWhiteSpace(c.ProductName) ? "FleetWarden" : c.ProductName.Trim());
            if (isNew)
            {
                lines.Children.Clear();
                lastLine = 0;
            }
            var techNew = false;
            foreach (var l in c.Lines ?? new ChatLine[0])
            {
                if (l.Id <= lastLine) continue;
                lastLine = l.Id;
                lines.Children.Add(Bubble(l));
                if (l.From == "tech") techNew = true;
            }
            compose.Visibility = c.Open ? Visibility.Visible : Visibility.Collapsed;
            if (!c.Open) ShowStatus(SsText.ChatEnded, false);
            else if (status.Text == SsText.ChatEnded) status.Visibility = Visibility.Collapsed;
            sendBtn.IsEnabled = !sending && c.Open && !string.IsNullOrWhiteSpace(input.Text);
            scroll.ScrollToEnd();
            return techNew || (isNew && c.Open);
        }

        FrameworkElement Bubble(ChatLine l)
        {
            var mine = l.From == "user";
            var sp = new StackPanel { Margin = new Thickness(mine ? 48 : 0, 0, mine ? 0 : 48, 10), HorizontalAlignment = mine ? HorizontalAlignment.Right : HorizontalAlignment.Left };
            var b = new Border { CornerRadius = mine ? new CornerRadius(12, 12, 4, 12) : new CornerRadius(12, 12, 12, 4), Padding = new Thickness(12, 8, 12, 8) };
            var t = new TextBlock { Text = l.Text ?? "", TextWrapping = TextWrapping.Wrap, LineHeight = 19 };
            if (mine)
            {
                b.SetResourceReference(Border.BackgroundProperty, "Accent");
                t.SetResourceReference(TextBlock.ForegroundProperty, "AccentFg");
            }
            else
            {
                b.SetResourceReference(Border.BackgroundProperty, "Surface");
                b.SetResourceReference(Border.BorderBrushProperty, "Line");
                b.BorderThickness = new Thickness(1);
            }
            b.Child = t;
            var meta = new TextBlock { Text = (mine ? SsText.ChatYou : l.Name) + " · " + Time(l.At), FontSize = 11, Margin = new Thickness(4, 0, 4, 3),
                HorizontalAlignment = mine ? HorizontalAlignment.Right : HorizontalAlignment.Left };
            meta.SetResourceReference(TextBlock.ForegroundProperty, "Muted");
            sp.Children.Add(meta);
            sp.Children.Add(b);
            return sp;
        }

        static string Time(string iso)
        {
            if (string.IsNullOrEmpty(iso) || !DateTime.TryParse(iso, CultureInfo.InvariantCulture, DateTimeStyles.AdjustToUniversal | DateTimeStyles.AssumeUniversal, out var t))
                return "";
            return t.ToLocalTime().ToString("HH:mm", CultureInfo.InvariantCulture);
        }

        async Task SendInput()
        {
            var text = (input.Text ?? "").Trim();
            if (text == "" || sending || State == null || !State.Open) return;
            sending = true;
            sendBtn.IsEnabled = false;
            SelfServiceReply r;
            try { r = await send(text); }
            catch (Exception) { r = new SelfServiceReply { Error = "offline" }; }
            sending = false;
            if (r == null || !string.IsNullOrEmpty(r.Error))
            {
                ShowStatus(!string.IsNullOrEmpty(r?.Message) ? r.Message : SsText.Error(r?.Error ?? "offline"), true);
                sendBtn.IsEnabled = !string.IsNullOrWhiteSpace(input.Text);
                return;
            }
            input.Text = "";
            status.Visibility = Visibility.Collapsed;
            if (r.Catalog?.Chat != null) Apply(r.Catalog.Chat);
            input.Focus();
        }

        void ShowStatus(string text, bool error)
        {
            status.Text = text;
            status.SetResourceReference(TextBlock.ForegroundProperty, error ? "Danger" : "Muted");
            status.Visibility = Visibility.Visible;
        }

        // Bring shows the window and draws attention without stealing the
        // keyboard from what the user is typing elsewhere.
        public void Bring()
        {
            if (!IsVisible)
            {
                ShowActivated = false;
                Show();
            }
            if (WindowState == WindowState.Minimized) WindowState = WindowState.Normal;
            scroll.ScrollToEnd();
        }

        public void CloseQuietly()
        {
            quitting = true;
            Close();
        }

        protected override void OnClosing(CancelEventArgs e)
        {
            if (!quitting)
            {
                e.Cancel = true;
                Hide();
            }
            base.OnClosing(e);
        }
    }
}
