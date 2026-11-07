using System;
using System.ComponentModel;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Markup;
using System.Windows.Media;

namespace FleetWarden.Tray
{
    // The bar at the top of the screen while a remote session runs. It stays
    // on top and cannot be closed, except with its button, which ends the
    // session.
    public sealed class RemoteBanner : Window
    {
        const string Layout = @"
<Border xmlns='http://schemas.microsoft.com/winfx/2006/xaml/presentation'
        xmlns:x='http://schemas.microsoft.com/winfx/2006/xaml'
        Margin='12' CornerRadius='10' Background='{DynamicResource Surface}' BorderBrush='{DynamicResource Accent}' BorderThickness='1.5'
        TextElement.FontFamily='Segoe UI Variable Text, Segoe UI' TextElement.Foreground='{DynamicResource Fg}'>
  <Border.Effect>
    <DropShadowEffect BlurRadius='20' ShadowDepth='4' Direction='270' Opacity='0.25' Color='#000000'/>
  </Border.Effect>
  <Border.Resources>
    <Style x:Key='Btn' TargetType='Button'>
      <Setter Property='Cursor' Value='Hand'/>
      <Setter Property='Height' Value='28'/>
      <Setter Property='Padding' Value='12,0'/>
      <Setter Property='FontSize' Value='12'/>
      <Setter Property='FontWeight' Value='SemiBold'/>
      <Setter Property='BorderThickness' Value='1'/>
      <Setter Property='FocusVisualStyle' Value='{x:Null}'/>
      <Setter Property='Template'>
        <Setter.Value>
          <ControlTemplate TargetType='Button'>
            <Border x:Name='Bd' CornerRadius='7' Background='{TemplateBinding Background}' BorderBrush='{TemplateBinding BorderBrush}'
                    BorderThickness='{TemplateBinding BorderThickness}' Padding='{TemplateBinding Padding}'>
              <ContentPresenter HorizontalAlignment='Center' VerticalAlignment='Center'/>
            </Border>
            <ControlTemplate.Triggers>
              <Trigger Property='IsMouseOver' Value='True'><Setter TargetName='Bd' Property='Opacity' Value='0.88'/></Trigger>
              <Trigger Property='IsPressed' Value='True'><Setter TargetName='Bd' Property='Opacity' Value='0.75'/></Trigger>
            </ControlTemplate.Triggers>
          </ControlTemplate>
        </Setter.Value>
      </Setter>
    </Style>
  </Border.Resources>
  <StackPanel Orientation='Horizontal' Margin='14,8,8,8'>
    <Ellipse Width='9' Height='9' Fill='#E5484D' VerticalAlignment='Center'/>
    <TextBlock x:Name='Text' Margin='10,0,14,0' VerticalAlignment='Center' FontSize='13' FontWeight='SemiBold'/>
    <TextBlock x:Name='Product' Margin='0,0,14,0' VerticalAlignment='Center' FontSize='12' Foreground='{DynamicResource Muted}'/>
    <Button x:Name='End' Style='{StaticResource Btn}' Background='{DynamicResource Accent}' BorderBrush='{DynamicResource Accent}' Foreground='{DynamicResource AccentFg}'/>
  </StackPanel>
</Border>";

        public string SessionId { get; }
        public Prompt Prompt { get; }
        bool closing;

        public RemoteBanner(string id, Prompt prompt, Action onEnd)
        {
            SessionId = id;
            Prompt = prompt;
            Title = Strings.Product(prompt);
            WindowStyle = WindowStyle.None;
            AllowsTransparency = true;
            Background = Brushes.Transparent;
            ResizeMode = ResizeMode.NoResize;
            Topmost = true;
            ShowInTaskbar = false;
            ShowActivated = false;
            SizeToContent = SizeToContent.WidthAndHeight;
            Theme.Apply(Resources, prompt.AccentColor);

            var root = (FrameworkElement)XamlReader.Parse(Layout);
            Content = root;
            ((TextBlock)root.FindName("Text")).Text = Strings.BannerText(prompt);
            ((TextBlock)root.FindName("Product")).Text = Strings.Product(prompt);
            var end = (Button)root.FindName("End");
            end.Content = Strings.EndSession;
            end.Click += (s, e) => { end.IsEnabled = false; onEnd?.Invoke(); };
            Loaded += (s, e) => Place();
            SizeChanged += (s, e) => { if (IsLoaded) Place(); };
        }

        void Place()
        {
            var area = SystemParameters.WorkArea;
            Left = area.Left + (area.Width - ActualWidth) / 2;
            Top = area.Top;
        }

        public void CloseQuietly()
        {
            closing = true;
            Close();
        }

        protected override void OnClosing(CancelEventArgs e)
        {
            if (!closing) e.Cancel = true;
            base.OnClosing(e);
        }
    }
}
